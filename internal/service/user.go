package service

import (
	"fmt"
	"github.com/jianfengye/collection"
	"github.com/jinzhu/copier"
	"github.com/towelong/lin-cms-go/internal/domain/dto"
	"github.com/towelong/lin-cms-go/internal/domain/model"
	"github.com/towelong/lin-cms-go/internal/domain/vo"
	"github.com/towelong/lin-cms-go/pkg"
	"github.com/towelong/lin-cms-go/pkg/response"
	"gorm.io/gorm"
)

type IUserService interface {
	GetUserById(id int) (model.User, error)
	GetUserByUsername(username string) (model.User, error)
	GetUserPageByGroupId(groupId int, page int, count int) (*vo.Page, error)
	IsAdmin(id int) (bool, error)
	VerifyUser(username, password string) (model.User, error)
	GetRootUserId() int
	ChangeUserPassword(id int, newPassword string) error
	DeleteUser(id int) error
	CreateUser(dto dto.RegisterDTO) error
	CreateUsernamePasswordIdentity(userId int, username, password string) error
}

type UserService struct {
	DB           *gorm.DB
	GroupService GroupService
}

func (u UserService) GetRootUserId() int {
	var (
		group     model.Group
		userGroup model.UserGroup
	)
	err := u.DB.Where("level = ?", Root).First(&group).Error
	if err != nil {
		return 0
	}
	err = u.DB.Where("group_id = ?", group.ID).First(&userGroup).Error
	if err != nil {
		return 0
	}
	return userGroup.UserID
}

func (u UserService) GetUserPageByGroupId(groupId int, page int, count int) (*vo.Page, error) {
	var (
		userGroups []model.UserGroup
		users      []model.User
		usersVo    []*vo.User
	)
	p := vo.NewPage(page, count)
	rootId := u.GetRootUserId()
	// groupId = 0 返回所有的分页用户
	if groupId == 0 {
		if err := u.DB.Where("user_id <> ?", rootId).Find(&userGroups).Error; err != nil {
			return p, err
		}
	} else {
		if err := u.DB.Where("user_id <> ? AND group_id = ?", rootId, groupId).Find(&userGroups).Error; err != nil {
			return p, err
		}
	}
	userGroupCollection := collection.NewObjCollection(userGroups)
	userIds, err := userGroupCollection.Pluck("UserID").ToInts()
	if err != nil {
		fmt.Println(err)
	}
	// 若非root用户数量为0，直接返回
	if len(userIds) == 0 {
		p.SetTotal(0)
		users = make([]model.User, 0)
		p.SetItems(users)
		return p, nil
	}
	db := u.DB.Limit(count).Offset(page*count).Find(&users, userIds)
	if db.Error != nil {
		return p, err
	}
	err = copier.Copy(&usersVo, &users)
	if err != nil {
		fmt.Println(err)
	}
	for _, user := range usersVo {
		groups, err := u.GroupService.GetUserGroupByUserId(user.ID)
		if err != nil {
			continue
		}
		var groupsVo []vo.Group
		err = copier.Copy(&groupsVo, &groups)
		if err != nil {
			fmt.Println(err)
		}
		user.Group = append(user.Group, groupsVo...)
	}
	p.SetItems(usersVo)
	p.SetTotal(int(db.RowsAffected))
	return p, nil
}

func (u UserService) GetUserById(id int) (model.User, error) {
	var user model.User
	res := u.DB.First(&user, "id = ?", id)
	if res.RowsAffected > 0 {
		return user, nil
	}
	return user, res.Error
}

func (u UserService) GetUserByUsername(username string) (model.User, error) {
	var user model.User
	err := u.DB.Where("username = ?", username).First(&user).Error
	return user, err
}

func (u UserService) GetUserByEmail(email string) (model.User, error) {
	var user model.User
	err := u.DB.Where("email = ?", email).First(&user).Error
	return user, err
}

func (u UserService) IsAdmin(id int) (bool, error) {
	// 先判断用户是否存在
	user, err := u.GetUserById(id)
	if err != nil {
		return false, err
	}
	// 查找root用户的分组id
	group, groupErr := u.GroupService.GetGroupByLevel(Root)
	if groupErr != nil {
		return false, groupErr
	}
	// 查询用户分组表中是否存在记录
	var userGroup model.UserGroup
	res := u.DB.Where("user_id = ? AND group_id = ?", user.ID, group.ID).First(&userGroup)
	if res.RowsAffected > 0 {
		return true, nil
	}
	return false, res.Error
}

func (u UserService) VerifyUser(username, password string) (model.User, error) {
	var (
		userIdentity model.UserIdentity
		user         model.User
	)
	db := u.DB.Where("identity_type = ? AND identifier = ?", UserPassword.String(), username).First(&userIdentity)
	if db.Error != nil {
		return user, response.NewResponse(10031)
	}
	verifyPsw := pkg.VerifyPsw(password, userIdentity.Credential)
	if verifyPsw {
		err := u.DB.Where("username = ?", username).First(&user).Error
		if err != nil {
			return user, response.NewResponse(10031)
		}
		return user, nil
	}
	return user, response.NewResponse(10032)
}

func (u UserService) ChangeUserPassword(id int, newPassword string) error {
	user, err := u.GetUserById(id)
	if err != nil {
		return response.NewResponse(10021)
	}
	var userIdentity model.UserIdentity
	db := u.DB.Where("user_id = ?", user.ID).First(&userIdentity)
	password := pkg.EncodePassword(newPassword)
	save := db.Debug().Model(&userIdentity).Update("credential", password)
	return save.Error
}

func (u UserService) DeleteUser(id int) error {
	user, err := u.GetUserById(id)
	if err != nil {
		return response.NewResponse(10021)
	}
	if u.GetRootUserId() == id {
		return response.NewResponse(10079)
	}
	// 1. 软删除user表中的数据
	u.DB.Delete(&user)
	// 2. 软删除user—identity表中的数据
	var userIdentity model.UserIdentity
	u.DB.Where("user_id = ?", user.ID).Delete(&userIdentity)
	// 3. 软删除user-group表中的数据
	var userGroup model.UserGroup
	update := u.DB.Where("user_id = ?", user.ID).Delete(&userGroup)
	return update.Error
}

func (u *UserService) CreateUser(dto dto.RegisterDTO) error {
	user, err := u.GetUserByUsername(dto.Username)
	// 若记录存在
	if user.ID > 0 {
		return response.NewResponse(10071)
	}
	if dto.Email != "" {
		userByEmail, _ := u.GetUserByEmail(dto.Email)
		// 若记录存在
		if userByEmail.ID > 0 {
			return response.NewResponse(10076)
		}
	}
	// 开启事务
	err = u.DB.Transaction(func(tx *gorm.DB) error {
		var user model.User
		copier.Copy(&user, &dto)
		if err := tx.Select("Username", "Email").Create(&user).Error; err != nil {
			return err
		}
		// 若指定了权限分组
		if dto.GroupIds != nil && len(dto.GroupIds) > 0 {
			if err := u.GroupService.CheckGroupsExist(dto.GroupIds); err != nil {
				return err
			}
			if err := u.GroupService.CheckGroupsValid(dto.GroupIds); err != nil {
				return err
			}
			var (
				userGroups = make([]model.UserGroup, 0)
				userGroup  model.UserGroup
			)
			for _, groupId := range dto.GroupIds {
				userGroups = append(userGroups, model.UserGroup{
					UserID:  user.ID,
					GroupID: groupId,
				})
			}
			if err := tx.Debug().Model(&userGroup).Create(userGroups).Error; err != nil {
				return err
			}
		} else {
			// 未指定分组则默认Guest
			guest, _ := u.GroupService.GetGroupByLevel(Guest)
			group := model.UserGroup{
				UserID:  user.ID,
				GroupID: guest.ID,
			}
			if err := tx.Create(&group).Error; err != nil {
				return err
			}
		}
		if err := u.CreateUsernamePasswordIdentity(user.ID, dto.Username, dto.Password); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return response.NewResponse(10200)
	}
	return nil
}

func (u *UserService) CreateUsernamePasswordIdentity(userId int, username, password string) error {
	userIdentity := model.UserIdentity{
		UserID:       userId,
		Identifier:   username,
		Credential:   pkg.EncodePassword(password),
		IdentityType: UserPassword.String(),
	}
	return u.DB.Select("UserID", "Identifier", "Credential", "IdentityType").Create(&userIdentity).Error
}