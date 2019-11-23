import * as React from 'react'
import {Link} from 'react-router-dom'
import {Layout, Icon, Menu} from 'antd'

import {MENUS} from '../../config/menu'
import StoreManager from '../../store/StoreManager'
import {BreadcrumbStatus} from '../../model/Breadcrumb'
import {autobind} from 'core-decorators'

const {Sider} = Layout
const Logo = require('../../assets/images/logo_title_dark.png')

interface SiderMenuProps {
}

interface SiderMenuStatus {
    currentBreadcrumbPath: Array<string>
}

export default class SiderMenu extends React.Component<SiderMenuProps, SiderMenuStatus> {
    breadcrumbStore: any
    flatMenu: Array<any>
    currentBreadcrumbPath: Array<any>

    constructor(props: SiderMenuProps) {
        super(props)
        this.breadcrumbStore = StoreManager.BreadcrumbStore
        this.flatMenu = this.getFlatMenu()
        this.currentBreadcrumbPath = []
        this.state = {
            currentBreadcrumbPath: []
        }
    }

    renderMenu(menus: Array<any>, parentPath?: string) {
        const IconTitle = (icon: string, title: string) => <span><Icon type={icon}/>{title}</span>

        return menus.map(menu => {
            const path = parentPath ? parentPath + menu.path : menu.path

            return menu.children
                ? (
                    <Menu.SubMenu key={menu.path} title={IconTitle(menu.icon, menu.title)}>
                        {this.renderMenu(menu.children, menu.path)}
                    </Menu.SubMenu>
                )
                : (
                    <Menu.Item key={path}>
                        <Link to={path}>{IconTitle(menu.icon, menu.title)}</Link>
                    </Menu.Item>
                )
        })
    }

    @autobind
    handleMenuClick(e: any) {
        /* e.keyPath为点击菜单所在的path以及父path组成的数组，父path在数组尾部*/
        this.currentBreadcrumbPath = e.keyPath.reverse()
        this.setBreadcrumbs()
    }

    @autobind
    setBreadcrumbs() {
        let breadcrumbs: Array<BreadcrumbStatus> = []
        this.currentBreadcrumbPath.map((path: string) => {
            this.flatMenu.map(item => {
                if (path == item.path) {
                    breadcrumbs.push(item)
                }
            })
        })
        this.breadcrumbStore.setBreadcrumbs(breadcrumbs)
    }

    @autobind
    getFlatMenu() {
        let flatMenu: Array<BreadcrumbStatus> = []
        MENUS.map(item => {
            if (item.children) {
                flatMenu.push({path: item.path, label: item.title})
                item.children.map(child => {
                    flatMenu.push({path: item.path + child.path, label: child.title})
                })
            } else {
                flatMenu.push({path: item.path, label: item.title})
            }
        })
        return flatMenu
    }

    @autobind
    initBreadcrumb() {
        const {location: {hash}} = window
        const path = hash.replace('#', '')
        let pathArr = path.split('/').slice(1)
        let result = ''
        let breadcrumbPathArray = pathArr.map((e, i) => {
            result += '/' + e
            return result
        })

        this.currentBreadcrumbPath = breadcrumbPathArray
        this.setBreadcrumbs()
    }

    componentDidMount(): void {
        this.initBreadcrumb()
    }

    render() {
        const {location: {hash}} = window
        const path = hash.replace('#', '')
        return (
            <Sider className="lindb-sider" collapsible={true} trigger={null}>
                {/* Logo */}
                <div className="lindb-sider__logo">
                    <Link to="/"><img src={Logo} alt="LinDB"/></Link>
                </div>

                {/* Menu */}
                <Menu
                    mode="inline"
                    theme="dark"
                    className="lindb-sider__menu"
                    defaultOpenKeys={['/monitoring', '/setting']}
                    selectedKeys={[path]}
                    onClick={this.handleMenuClick}
                >
                    {this.renderMenu(MENUS)}
                </Menu>
            </Sider>
        )
    }
}